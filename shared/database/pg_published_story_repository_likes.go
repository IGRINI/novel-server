package database

import (
	"context"
	"fmt"
	"novel-server/shared/models"

	"github.com/google/uuid"
	// "github.com/jackc/pgx/v5/pgxpool" // No longer needed here
	"go.uber.org/zap"
)

// Константы для операций с лайками
const (
	// Query to check if a like exists
	checkLikeExistsQuery = `SELECT EXISTS (SELECT 1 FROM story_likes WHERE user_id = $1 AND published_story_id = $2)`

	// Query to insert a like, ignoring conflicts (if like already exists)
	insertLikeQuery = `INSERT INTO story_likes (user_id, published_story_id, created_at) VALUES ($1, $2, NOW()) ON CONFLICT (user_id, published_story_id) DO NOTHING`

	// Query to delete a like
	deleteLikeQuery = `DELETE FROM story_likes WHERE user_id = $1 AND published_story_id = $2`

	// Query to increment the likes count on the published_stories table
	incrementLikesCountQuery = `UPDATE published_stories SET likes_count = likes_count + 1, updated_at = NOW() WHERE id = $1`

	// Query to decrement the likes count, ensuring it doesn't go below zero
	decrementLikesCountQuery = `UPDATE published_stories SET likes_count = GREATEST(0, likes_count - 1), updated_at = NOW() WHERE id = $1`

	// Query to update the like count directly (maybe less safe than increment/decrement?)
	updateLikeCountQuery = `UPDATE published_stories SET likes_count = $1, updated_at = NOW() WHERE id = $2`
)

// MarkStoryAsLiked отмечает историю как лайкнутую пользователем.
// ВНИМАНИЕ: Этот метод больше НЕ управляет транзакцией. Вызывающий код ДОЛЖЕН обернуть его в транзакцию.
func (r *pgPublishedStoryRepository) MarkStoryAsLiked(ctx context.Context, storyID uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Attempting to mark story as liked (transaction managed externally)", logFields...)

	// 1. Insert like record (ignore conflict)
	// Используем r.db напрямую, так как транзакция управляется извне.
	result, err := r.db.Exec(ctx, insertLikeQuery, userID, storyID)
	if err != nil {
		r.logger.Error("Failed to insert into story_likes", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка добавления лайка в story_likes: %w", err)
	}

	// 2. If inserted (RowsAffected > 0), increment counter
	if result.RowsAffected() > 0 {
		incrementResult, err := r.db.Exec(ctx, incrementLikesCountQuery, storyID)
		if err != nil {
			r.logger.Error("Failed to increment likes count after inserting like", append(logFields, zap.Error(err))...)
			// Не делаем rollback, так как транзакция внешняя
			return fmt.Errorf("ошибка инкремента счетчика лайков: %w", err)
		}
		if incrementResult.RowsAffected() == 0 {
			r.logger.Error("Story not found for incrementing likes count after inserting like record", logFields...)
			// Не делаем rollback
			return models.ErrNotFound // Story disappeared mid-operation?
		}
		r.logger.Debug("Likes count incremented", logFields...)
	} else {
		r.logger.Debug("Like record already existed, likes count not incremented", logFields...)
	}

	// Коммит или откат выполняется вызывающим кодом.
	r.logger.Info("MarkStoryAsLiked operation completed within external transaction context", logFields...)
	return nil
}

// MarkStoryAsUnliked отмечает историю как не лайкнутую пользователем.
// ВНИМАНИЕ: Этот метод больше НЕ управляет транзакцией. Вызывающий код ДОЛЖЕН обернуть его в транзакцию.
func (r *pgPublishedStoryRepository) MarkStoryAsUnliked(ctx context.Context, storyID uuid.UUID, userID uuid.UUID) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.String("userID", userID.String())}
	r.logger.Debug("Attempting to mark story as unliked (transaction managed externally)", logFields...)

	// 1. Delete like record
	result, err := r.db.Exec(ctx, deleteLikeQuery, userID, storyID)
	if err != nil {
		r.logger.Error("Failed to delete from story_likes", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка удаления лайка из story_likes: %w", err)
	}

	// 2. If deleted (RowsAffected > 0), decrement counter
	if result.RowsAffected() > 0 {
		decrementResult, err := r.db.Exec(ctx, decrementLikesCountQuery, storyID)
		if err != nil {
			r.logger.Error("Failed to decrement likes count after deleting like", append(logFields, zap.Error(err))...)
			return fmt.Errorf("ошибка декремента счетчика лайков: %w", err)
		}
		if decrementResult.RowsAffected() == 0 {
			r.logger.Error("Story not found for decrementing likes count after deleting like record", logFields...)
			return models.ErrNotFound // Story disappeared mid-operation?
		}
		r.logger.Debug("Likes count decremented", logFields...)
	} else {
		r.logger.Debug("Like record did not exist, likes count not decremented", logFields...)
	}

	// Коммит или откат выполняется вызывающим кодом.
	r.logger.Info("MarkStoryAsUnliked operation completed within external transaction context", logFields...)
	return nil
}

// UpdateLikeCount обновляет счетчик лайков для истории.
// Примечание: Обычно безопаснее использовать инкремент/декремент.
// Этот метод может быть полезен для синхронизации, если счетчик вычисляется отдельно.
func (r *pgPublishedStoryRepository) UpdateLikeCount(ctx context.Context, storyID uuid.UUID, count int64) error {
	logFields := []zap.Field{zap.String("storyID", storyID.String()), zap.Int64("newCount", count)}
	r.logger.Debug("Updating like count directly", logFields...)

	commandTag, err := r.db.Exec(ctx, updateLikeCountQuery, count, storyID)
	if err != nil {
		r.logger.Error("Failed to update like count", append(logFields, zap.Error(err))...)
		return fmt.Errorf("ошибка обновления счетчика лайков для истории %s: %w", storyID, err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("No rows affected when updating like count (story not found?)", logFields...)
		return models.ErrNotFound // Story not found
	}

	r.logger.Info("Like count updated successfully", logFields...)
	return nil
}

// CheckLike проверяет, лайкнул ли пользователь историю.
func (r *pgPublishedStoryRepository) CheckLike(ctx context.Context, userID, storyID uuid.UUID) (bool, error) {
	logFields := []zap.Field{zap.String("userID", userID.String()), zap.String("storyID", storyID.String())}
	r.logger.Debug("Checking like status", logFields...)

	var exists bool
	err := r.db.QueryRow(ctx, checkLikeExistsQuery, userID, storyID).Scan(&exists)
	if err != nil {
		// pgx.ErrNoRows should not happen with EXISTS, but handle defensively
		r.logger.Error("Error checking like status from DB", append(logFields, zap.Error(err))...)
		return false, fmt.Errorf("ошибка проверки лайка в БД: %w", err)
	}

	r.logger.Debug("Like status checked", append(logFields, zap.Bool("exists", exists))...)
	return exists, nil
}
