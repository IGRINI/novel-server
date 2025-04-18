package database

import (
	"context"
	"errors"
	"fmt"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

// pgLikeRepository реализует интерфейс LikeRepository для PostgreSQL.
type pgLikeRepository struct {
	db     interfaces.DBTX
	logger *zap.Logger
}

// Compile-time check
var _ interfaces.LikeRepository = (*pgLikeRepository)(nil)

// NewPgLikeRepository создает новый экземпляр репозитория лайков.
func NewPgLikeRepository(db interfaces.DBTX, logger *zap.Logger) interfaces.LikeRepository {
	return &pgLikeRepository{
		db:     db,
		logger: logger.Named("PgLikeRepo"),
	}
}

// AddLike добавляет запись о лайке.
func (r *pgLikeRepository) AddLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	query := `INSERT INTO story_likes (user_id, published_story_id) VALUES ($1, $2)`
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Adding like record", logFields...)

	_, err := r.db.Exec(ctx, query, userID, publishedStoryID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation (означает, что лайк уже существует)
				r.logger.Warn("Like already exists (unique constraint violation)", logFields...)
				return interfaces.ErrLikeAlreadyExists
			case "23503": // foreign_key_violation (означает, что published_story_id не найден)
				r.logger.Warn("Published story not found (foreign key violation)", logFields...)
				return models.ErrNotFound // Возвращаем стандартную ошибку
			}
		}
		// Другая ошибка БД
		r.logger.Error("Failed to add like record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to add like: %w", err)
	}

	r.logger.Info("Like record added successfully", logFields...)
	return nil
}

// RemoveLike удаляет запись о лайке.
func (r *pgLikeRepository) RemoveLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) error {
	query := `DELETE FROM story_likes WHERE user_id = $1 AND published_story_id = $2`
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Removing like record", logFields...)

	commandTag, err := r.db.Exec(ctx, query, userID, publishedStoryID)
	if err != nil {
		r.logger.Error("Failed to remove like record", append(logFields, zap.Error(err))...)
		return fmt.Errorf("failed to remove like: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		r.logger.Warn("Like not found to remove", logFields...)
		return interfaces.ErrLikeNotFound // Лайка не было
	}

	r.logger.Info("Like record removed successfully", logFields...)
	return nil
}

// CheckLike проверяет, лайкнул ли пользователь историю.
func (r *pgLikeRepository) CheckLike(ctx context.Context, userID uuid.UUID, publishedStoryID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS (SELECT 1 FROM story_likes WHERE user_id = $1 AND published_story_id = $2)`
	var exists bool
	logFields := []zap.Field{
		zap.String("userID", userID.String()),
		zap.String("publishedStoryID", publishedStoryID.String()),
	}
	r.logger.Debug("Checking if like exists", logFields...)

	err := r.db.QueryRow(ctx, query, userID, publishedStoryID).Scan(&exists)
	if err != nil {
		r.logger.Error("Failed to check like existence", append(logFields, zap.Error(err))...)
		return false, fmt.Errorf("failed to check like existence: %w", err)
	}

	r.logger.Debug("Like existence check completed", append(logFields, zap.Bool("exists", exists))...)
	return exists, nil
}

// CountLikes возвращает общее количество лайков для истории.
func (r *pgLikeRepository) CountLikes(ctx context.Context, publishedStoryID uuid.UUID) (int64, error) {
	query := `SELECT COUNT(*) FROM story_likes WHERE published_story_id = $1`
	var count int64
	logFields := []zap.Field{zap.String("publishedStoryID", publishedStoryID.String())}
	r.logger.Debug("Counting likes for story", logFields...)

	err := r.db.QueryRow(ctx, query, publishedStoryID).Scan(&count)
	if err != nil {
		// Если история не найдена, COUNT вернет 0, ошибки не будет (если FK не строгий или история удалена каскадно)
		// Поэтому проверяем на pgx.ErrNoRows здесь не нужно.
		r.logger.Error("Failed to count likes for story", append(logFields, zap.Error(err))...)
		return 0, fmt.Errorf("failed to count likes: %w", err)
	}

	r.logger.Debug("Likes counted successfully", append(logFields, zap.Int64("count", count))...)
	return count, nil
}
