package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// Константы для прочих операций
const (
	deleteStoryLikesQuery     = `DELETE FROM story_likes WHERE published_story_id = $1`
	deletePlayerProgressQuery = `DELETE FROM player_progress WHERE published_story_id = $1`
	deleteStoryScenesQuery    = `DELETE FROM story_scenes WHERE published_story_id = $1`
	deletePublishedStoryQuery = `DELETE FROM published_stories WHERE id = $1`
	incrementViewCountQuery   = `UPDATE published_stories SET view_count = view_count + 1, updated_at = NOW() WHERE id = $1`
	checkStoryExistsQuery     = `SELECT user_id FROM published_stories WHERE id = $1`
)

// Delete удаляет опубликованную историю и все связанные с ней данные.
// Требует userID для проверки владения.
// ВНИМАНИЕ: Этот метод больше НЕ управляет транзакцией. Вызывающий код ДОЛЖЕН обернуть его в транзакцию.
func (r *pgPublishedStoryRepository) Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{
		zap.String("publishedStoryID", id.String()),
		zap.String("userID", userID.String()),
	}
	r.logger.Info("Attempting to delete published story and related data (transaction managed externally)", logFields...)

	// Транзакция управляется извне, используем r.db напрямую

	// 1. Check ownership
	var ownerID uuid.UUID
	err := r.db.QueryRow(ctx, checkStoryExistsQuery, id).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			r.logger.Warn("Published story not found for deletion", logFields...)
			return models.ErrNotFound
		}
		r.logger.Error("Failed to check story ownership before deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка проверки владения историей %s: %w", id, err)
	}
	if ownerID != userID {
		r.logger.Warn("User does not own the story attempted for deletion", logFields...)
		return models.ErrForbidden
	}

	// 2. Delete related data (Order: likes -> progress -> states -> scenes -> story)
	// Используем r.db для всех операций

	// 2a. Delete likes
	if _, err := r.db.Exec(ctx, deleteStoryLikesQuery, id); err != nil {
		r.logger.Error("Failed to delete story_likes during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления лайков для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related likes", logFields...)

	// 2b. Delete player progress
	if _, err := r.db.Exec(ctx, deletePlayerProgressQuery, id); err != nil {
		r.logger.Error("Failed to delete player_progress during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления прогресса игроков для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related player progress", logFields...)

	// 2c. Delete player game states (assuming relation exists)
	// TODO: Add query and execution for deleting player_game_states if necessary
	// deleteGameStatesQuery := `DELETE FROM player_game_states WHERE published_story_id = $1`
	// if _, err := r.db.Exec(ctx, deleteGameStatesQuery, id); err != nil { ... }

	// 2d. Delete story scenes
	if _, err := r.db.Exec(ctx, deleteStoryScenesQuery, id); err != nil {
		r.logger.Error("Failed to delete story_scenes during story deletion", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления сцен для истории %s: %w", id, err)
	}
	r.logger.Debug("Deleted related scenes", logFields...)

	// 3. Delete the story itself
	commandTag, err := r.db.Exec(ctx, deletePublishedStoryQuery, id)
	if err != nil {
		r.logger.Error("Failed to delete published_stories record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления основной записи истории %s: %w", id, err)
	}
	if commandTag.RowsAffected() == 0 {
		// Should not happen as we checked existence
		r.logger.Error("Published story disappeared during deletion operation", logFields...)
		return models.ErrNotFound
	}
	r.logger.Debug("Deleted published_stories record", logFields...)

	// Коммит или откат выполняется вызывающим кодом.
	r.logger.Info("Published story and related data delete operations completed within external transaction context", logFields...)
	return nil
}

// IncrementViewCount увеличивает счетчик просмотров для истории.
func (r *pgPublishedStoryRepository) IncrementViewCount(ctx context.Context, storyID uuid.UUID) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String())}
	r.logger.Debug("Incrementing view count", logFields...)

	commandTag, err := r.db.Exec(ctx, incrementViewCountQuery, storyID)
	if err != nil {
		// Log the error but don't necessarily return a fatal error to the user,
		// as failing to increment view count might not be critical.
		r.logger.Error("Failed to increment view count", append(logFields, zap.Error(err))...)
		// Depending on requirements, might return nil or the error
		return fmt.Errorf("ошибка инкремента счетчика просмотров для истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when incrementing view count (story not found?)", logFields...)
		// Story not found is a valid case where count doesn't increment.
		// Depending on requirements, might return ErrNotFound or nil.
		return models.ErrNotFound
	}

	r.logger.Debug("View count incremented successfully", logFields...)
	return nil
}
